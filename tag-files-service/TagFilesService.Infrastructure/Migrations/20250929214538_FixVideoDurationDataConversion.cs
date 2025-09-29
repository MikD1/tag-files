using Microsoft.EntityFrameworkCore.Migrations;

#nullable disable

namespace TagFilesService.Infrastructure.Migrations
{
    /// <inheritdoc />
    public partial class FixVideoDurationDataConversion : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.Sql(@"
                UPDATE LibraryItems
                SET VideoDuration =
                    (
                        /* whole seconds -> ticks */
                        (
                            CAST(SUBSTR(VideoDuration,1,2) AS INTEGER) * 3600 +
                            CAST(SUBSTR(VideoDuration,4,2) AS INTEGER) * 60  +
                            CAST(SUBSTR(VideoDuration,7,2) AS INTEGER)
                        ) * 10000000
                    )
                    +
                    /* fractional part -> ticks (pad/truncate to 7 digits) */
                    CASE
                        WHEN INSTR(VideoDuration, '.') > 0 THEN
                            CAST(
                                SUBSTR(
                                    (SUBSTR(VideoDuration, INSTR(VideoDuration, '.') + 1) || '0000000'),
                                    1, 7
                                ) AS INTEGER
                            )
                        ELSE 0
                    END
                WHERE typeof(VideoDuration) = 'text'
                      AND VideoDuration IS NOT NULL;
            ");
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
        }
    }
}
