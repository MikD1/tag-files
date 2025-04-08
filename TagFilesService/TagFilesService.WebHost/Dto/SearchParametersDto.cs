namespace TagFilesService.WebHost.Dto;

public record SearchParametersDto(
    string TagQuery,
    int PageIndex,
    int PageSize);