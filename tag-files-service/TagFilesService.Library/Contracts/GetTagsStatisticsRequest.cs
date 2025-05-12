using MediatR;

namespace TagFilesService.Library.Contracts;

public record GetTagsStatisticsRequest : IRequest<List<TagStatisticsDto>>;